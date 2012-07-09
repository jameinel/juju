package mstate

import (
	"fmt"
	"labix.org/v2/mgo/bson"
)

// Machine represents the state of a machine.
type Machine struct {
	st *State
	id int
}

// machineDoc represents the internal state of a machine in MongoDB.
type machineDoc struct {
	Id         int `bson:"_id"`
	InstanceId string
	Life       Life
}

// Id returns the machine id.
func (m *Machine) Id() int {
	return m.id
}

// InstanceId returns the provider specific machine id for this machine.
func (m *Machine) InstanceId() (string, error) {
	mdoc := &machineDoc{}
	sel := bson.D{
		{"_id", m.id},
		{"life", Alive},
	}
	err := m.st.machines.Find(sel).One(mdoc)
	if err != nil {
		return "", fmt.Errorf("can't get instance id of machine %s: %v", m, err)
	}
	return mdoc.InstanceId, nil
}

// Units returns all the units that have been assigned to the machine.
func (m *Machine) Units() (units []*Unit, err error) {
	defer errorContextf(&err, "can't get units assigned to machine %s", m)
	pudocs := []unitDoc{}
	err = m.st.units.Find(bson.D{{"machineid", m.id}}).All(&pudocs)
	if err != nil {
		return nil, err
	}
	for _, pudoc := range pudocs {
		docs := []unitDoc{}
		sel := bson.D{{"principal", pudoc.Name}, {"life", Alive}}
		err = m.st.units.Find(sel).All(&docs)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			units = append(units, newUnit(m.st, &doc))
		}
	}
	return units, nil
}

// SetInstanceId sets the provider specific machine id for this machine.
func (m *Machine) SetInstanceId(id string) error {
	change := bson.D{{"$set", bson.D{{"instanceid", id}}}}
	err := m.st.machines.Update(bson.D{{"_id", m.id}}, change)
	if err != nil {
		return fmt.Errorf("can't set instance id of machine %s: %v", m, err)
	}
	return nil
}

func machineNameFromId(id int) string {
	return fmt.Sprintf("machine-%010d", id)
}

// String returns a unique description of this machine.
func (m *Machine) String() string {
	return machineNameFromId(m.id)
}

func (m *machineDoc) String() string {
	return machineNameFromId(m.Id)
}
